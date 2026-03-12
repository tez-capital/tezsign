# Add our custom function to the end of the image generation pipeline
IMAGE_POSTPROCESS_COMMAND += "extract_final_image;"

extract_final_image() {
    # Create a 'release' directory next to the kas.yml file
    mkdir -p ${TOPDIR}/../release
    
    # Copy the WIC file and rename it to gadget-os.img
    if [ -e ${IMGDEPLOYDIR}/${IMAGE_LINK_NAME}.wic ]; then
        cp ${IMGDEPLOYDIR}/${IMAGE_LINK_NAME}.wic ${TOPDIR}/../release/gadget-os.img
        echo "=================================================================="
        echo "SUCCESS: Your final flashable image is at release/gadget-os.img"
        echo "=================================================================="
    fi
}